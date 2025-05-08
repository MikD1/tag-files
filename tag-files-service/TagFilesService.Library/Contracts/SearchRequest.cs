using MediatR;
using TagFilesService.Model;

namespace TagFilesService.Library.Contracts;

public record SearchRequest(
    string? TagQuery,
    FileType? ItemType,
    int PageIndex,
    int PageSize)
    : IRequest<PaginatedList<LibraryItemDto>>;