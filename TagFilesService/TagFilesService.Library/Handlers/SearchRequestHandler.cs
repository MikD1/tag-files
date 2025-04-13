using MediatR;
using TagFilesService.Library.Contracts;

namespace TagFilesService.Library.Handlers;

internal class SearchRequestHandler : IRequestHandler<SearchRequest>
{
    public Task Handle(SearchRequest request, CancellationToken cancellationToken)
    {
        throw new NotImplementedException();
    }
}