using Microsoft.EntityFrameworkCore;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class PaginatedList<T> : IPaginatedList<T>
{
    public static PaginatedList<T> Create(IQueryable<T> source, int pageIndex, int pageSize)
    {
        int count = source.Count();
        IQueryable<T> query = MakeQuery(source, pageIndex, pageSize);
        List<T> items = query.ToList();
        return new(items, count, pageIndex, pageSize);
    }

    public static async Task<PaginatedList<T>> CreateAsync(IQueryable<T> source, int pageIndex, int pageSize)
    {
        int count = await source.CountAsync();
        IQueryable<T> query = MakeQuery(source, pageIndex, pageSize);
        List<T> items = await query.ToListAsync();
        return new(items, count, pageIndex, pageSize);
    }

    public List<T> Items { get; }

    public int TotalItems { get; }

    public int PageIndex { get; }

    public int TotalPages { get; }

    private static IQueryable<T> MakeQuery(IQueryable<T> source, int pageIndex, int pageSize)
    {
        return source.Skip((pageIndex - 1) * pageSize).Take(pageSize);
    }

    private PaginatedList(List<T> items, int totalItems, int pageIndex, int pageSize)
    {
        Items = items;
        TotalItems = totalItems;
        PageIndex = pageIndex;
        TotalPages = (int)Math.Ceiling(totalItems / (double)pageSize);
    }
}